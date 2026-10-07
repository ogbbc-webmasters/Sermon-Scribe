port module Main exposing (main)

import Api
import Browser
import Browser.Navigation as Navigation
import Dict
import Http
import Json.Decode as Decode
import Set
import Task
import Time
import Types exposing (Model, Msg(..), SermonList(..), UploadState(..))
import Url exposing (Url)
import Url.Parser as Parser exposing ((</>), s, string)
import View


port pipelineEvents : (Decode.Value -> msg) -> Sub msg


port copyTranscript : String -> Cmd msg


port transcriptCopied : (Bool -> msg) -> Sub msg


port scrollTranscriptMatch : Int -> Cmd msg


main : Program () Model Msg
main =
    Browser.application
        { init = init
        , update = update
        , view = \model -> { title = "Sermon Scribe", body = [ View.view model ] }
        , subscriptions = subscriptions
        , onUrlRequest = UrlRequested
        , onUrlChange = UrlChanged
        }


init : () -> Url -> Navigation.Key -> ( Model, Cmd Msg )
init _ url key =
    ( { sermons = Loading
      , selectedSermon = sermonIdFromUrl url
      , navigationKey = key
      , transcriptSearch = ""
      , transcriptMatch = 0
      , transcriptCopyStatus = Nothing
      , hasPipelineSnapshot = False
      , upload = Idle
      , confirmingDelete = Nothing
      , deleting = Set.empty
      , deletedSermons = Set.empty
      , deleteError = Nothing
      , retrying = Set.empty
      , regenerating = Dict.empty
      , retryError = Nothing
      , rerunning = Set.empty
      , reviewingNormalization = Set.empty
      , normalizationError = Nothing
      , zone = Time.utc
      }
    , Cmd.batch [ Api.fetchSermons GotSermons, Task.perform GotZone Time.here ]
    )



-- UPDATE


sermonIdFromUrl : Url -> Maybe String
sermonIdFromUrl url =
    Parser.parse (s "sermons" </> string) url


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        NoOp ->
            ( model, Cmd.none )

        UrlRequested request ->
            case request of
                Browser.Internal url ->
                    ( model, Navigation.pushUrl model.navigationKey (Url.toString url) )

                Browser.External url ->
                    ( model, Navigation.load url )

        UrlChanged url ->
            ( { model
                | selectedSermon = sermonIdFromUrl url
                , transcriptSearch = ""
                , transcriptMatch = 0
                , transcriptCopyStatus = Nothing
                , confirmingDelete = Nothing
                , retryError = Nothing
                , deleteError = Nothing
              }
            , Cmd.none
            )

        GotZone zone ->
            ( { model | zone = zone }, Cmd.none )

        GotSermons (Ok sermons) ->
            if model.hasPipelineSnapshot then
                ( model, Cmd.none )

            else
                ( { model | sermons = mergeFetchedSermons model.deletedSermons sermons model.sermons }
                , Cmd.none
                )

        GotSermons (Err _) ->
            if model.hasPipelineSnapshot then
                ( model, Cmd.none )

            else
                ( { model | sermons = LoadFailed }, Cmd.none )

        PipelineEventReceived value ->
            case Decode.decodeValue Api.pipelineEventDecoder value of
                Ok (Api.PipelineSnapshot sermons) ->
                    let
                        visible =
                            List.filter (\sermon -> not (Set.member sermon.id model.deletedSermons)) sermons

                        updated =
                            { model
                                | sermons =
                                    Loaded visible
                                , hasPipelineSnapshot = True
                            }
                    in
                    ( updated, Cmd.none )

                Ok (Api.PipelineUpdate sermon) ->
                    if Set.member sermon.id model.deletedSermons then
                        ( model, Cmd.none )

                    else
                        ( { model | sermons = upsertSermon sermon model.sermons }, Cmd.none )

                Ok (Api.PipelineDeleted id) ->
                    ( { model
                        | deleting = Set.remove id model.deleting
                        , deletedSermons = Set.insert id model.deletedSermons
                        , sermons = removeSermon id model.sermons
                      }
                    , Cmd.none
                    )

                Err _ ->
                    ( model, Cmd.none )

        FilePicked file ->
            ( { model | upload = Uploading 0 }
            , Api.uploadSermon UploadFinished file
            )

        UploadProgress progress ->
            case progress of
                Http.Sending sent ->
                    ( { model | upload = Uploading (Http.fractionSent sent) }
                    , Cmd.none
                    )

                Http.Receiving _ ->
                    ( model, Cmd.none )

        UploadFinished (Ok sermon) ->
            ( { model
                | upload = Idle
                , deleteError = Nothing
                , sermons = insertSermonIfMissing sermon model.sermons
              }
            , Cmd.none
            )

        UploadFinished (Err err) ->
            ( { model | upload = UploadFailed (uploadErrorMessage err) }
            , Cmd.none
            )

        RetryProcessing sermon part ->
            ( { model
                | retrying = Set.insert sermon.id model.retrying
                , regenerating = Dict.insert sermon.id part model.regenerating
                , retryError = Nothing
              }
            , Api.retryProcessing (RetryFinished sermon) sermon.id part
            )

        RetryFinished original (Ok sermon) ->
            ( { model
                | retrying = Set.remove original.id model.retrying
                , regenerating = Dict.remove original.id model.regenerating
                , retryError = Nothing
                , sermons =
                    if Set.member original.id model.deletedSermons then
                        model.sermons

                    else
                        replaceSermonIfUnchanged original sermon model.sermons
              }
            , Cmd.none
            )

        RetryFinished original (Err _) ->
            ( { model
                | retrying = Set.remove original.id model.retrying
                , regenerating = Dict.remove original.id model.regenerating
                , retryError = Just "Could not regenerate. Please try again."
              }
            , Cmd.none
            )

        RerunNormalization sermon adjustment ->
            ( { model
                | rerunning = Set.insert sermon.id model.rerunning
                , normalizationError = Nothing
              }
            , Api.rerunNormalization (RerunNormalizationFinished sermon) sermon.id adjustment
            )

        RerunNormalizationFinished original (Ok sermon) ->
            ( { model
                | rerunning = Set.remove original.id model.rerunning
                , normalizationError = Nothing
                , sermons =
                    if Set.member original.id model.deletedSermons then
                        model.sermons

                    else
                        replaceSermonIfUnchanged original sermon model.sermons
              }
            , Cmd.none
            )

        RerunNormalizationFinished original (Err _) ->
            ( { model
                | rerunning = Set.remove original.id model.rerunning
                , normalizationError = Just "Could not adjust the recording. Please try again."
              }
            , Cmd.none
            )

        ReviewNormalization sermon ->
            ( { model
                | reviewingNormalization = Set.insert sermon.id model.reviewingNormalization
                , normalizationError = Nothing
              }
            , Api.reviewNormalization (NormalizationReviewed sermon) sermon.id
            )

        NormalizationReviewed _ (Ok sermon) ->
            ( { model
                | sermons =
                    if Set.member sermon.id model.deletedSermons then
                        model.sermons

                    else
                        upsertSermon sermon model.sermons
                , reviewingNormalization = Set.remove sermon.id model.reviewingNormalization
              }
            , Cmd.none
            )

        NormalizationReviewed original (Err _) ->
            ( { model
                | reviewingNormalization = Set.remove original.id model.reviewingNormalization
                , normalizationError = Just "Could not save the normalization review. Please try again."
              }
            , Cmd.none
            )

        OpenSermon sermon ->
            ( model, Navigation.pushUrl model.navigationKey ("/sermons/" ++ Url.percentEncode sermon.id) )

        SearchTranscript query ->
            ( { model | transcriptSearch = query, transcriptMatch = 0 }, scrollTranscriptMatch 0 )

        SelectTranscriptMatch index ->
            ( { model | transcriptMatch = index }, scrollTranscriptMatch index )

        CopyTranscript transcript ->
            ( { model | transcriptCopyStatus = Nothing }, copyTranscript transcript )

        TranscriptCopied succeeded ->
            ( { model | transcriptCopyStatus = Just succeeded }, Cmd.none )

        AskDelete sermon ->
            ( { model | confirmingDelete = Just sermon }, Cmd.none )

        CancelDelete ->
            ( { model | confirmingDelete = Nothing }, Cmd.none )

        ConfirmDelete sermon ->
            ( { model
                | confirmingDelete = Nothing
                , deleting = Set.insert sermon.id model.deleting
                , deleteError = Nothing
              }
            , Api.deleteSermon (DeleteFinished sermon.id) sermon.id
            )

        DeleteFinished id (Ok ()) ->
            ( { model
                | deleting = Set.remove id model.deleting
                , deletedSermons = Set.insert id model.deletedSermons
                , deleteError = Nothing
                , sermons = removeSermon id model.sermons
              }
            , Cmd.none
            )

        DeleteFinished id (Err _) ->
            ( { model
                | deleting = Set.remove id model.deleting
                , deleteError = Just "Could not delete. Please try again."
              }
            , Cmd.none
            )


subscriptions : Model -> Sub Msg
subscriptions model =
    Sub.batch
        [ pipelineEvents PipelineEventReceived
        , transcriptCopied TranscriptCopied
        , case model.upload of
            Uploading _ ->
                Http.track Api.uploadTracker UploadProgress

            _ ->
                Sub.none
        ]


mergeFetchedSermons : Set.Set String -> List Api.Sermon -> SermonList -> SermonList
mergeFetchedSermons deleted fetched current =
    let
        visibleFetched =
            List.filter (\sermon -> not (Set.member sermon.id deleted)) fetched
    in
    case current of
        Loaded currentSermons ->
            List.foldl upsertSermon (Loaded visibleFetched) currentSermons

        _ ->
            Loaded visibleFetched


upsertSermon : Api.Sermon -> SermonList -> SermonList
upsertSermon sermon sermonList =
    case sermonList of
        Loaded sermons ->
            if List.any (\existing -> existing.id == sermon.id) sermons then
                Loaded
                    (List.map
                        (\existing ->
                            if existing.id == sermon.id then
                                sermon

                            else
                                existing
                        )
                        sermons
                    )

            else
                Loaded (sermon :: sermons)

        _ ->
            Loaded [ sermon ]


insertSermonIfMissing : Api.Sermon -> SermonList -> SermonList
insertSermonIfMissing sermon sermonList =
    case sermonList of
        Loaded sermons ->
            if List.any (\existing -> existing.id == sermon.id) sermons then
                sermonList

            else
                Loaded (sermon :: sermons)

        _ ->
            Loaded [ sermon ]


replaceSermonIfUnchanged : Api.Sermon -> Api.Sermon -> SermonList -> SermonList
replaceSermonIfUnchanged original replacement sermonList =
    case sermonList of
        Loaded sermons ->
            Loaded
                (List.map
                    (\existing ->
                        if existing == original then
                            replacement

                        else
                            existing
                    )
                    sermons
                )

        _ ->
            sermonList


removeSermon : String -> SermonList -> SermonList
removeSermon id sermonList =
    case sermonList of
        Loaded sermons ->
            Loaded (List.filter (\sermon -> sermon.id /= id) sermons)

        _ ->
            sermonList


uploadErrorMessage : Http.Error -> String
uploadErrorMessage err =
    case err of
        Http.BadStatus 413 ->
            "That file is too large. The limit is 2 GB."

        Http.NetworkError ->
            "The connection was interrupted. Please try again."

        Http.Timeout ->
            "The upload took too long. Please try again."

        _ ->
            "Something went wrong with the upload. Please try again."
