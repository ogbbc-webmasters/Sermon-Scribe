port module Main exposing (main)

import Api
import Browser
import Browser.Navigation as Navigation
import Dict
import Editing
import File.Select
import Http
import Json.Decode as Decode
import Json.Encode as Encode
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


port editingAudio : Encode.Value -> Cmd msg


port editingAudioStatus : (String -> msg) -> Sub msg


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
      , scriptureDrafts = Dict.empty
      , scriptureSaving = Set.empty
      , scriptureSaveErrors = Set.empty
      , zone = Time.utc
      , editor = Editing.init
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

        EditingMsg editorMsg ->
            let
                ( editor, cmd, audioEffect ) =
                    Editing.update editorMsg model.editor
            in
            ( { model | editor = editor }
            , Cmd.batch
                [ Cmd.map EditingMsg cmd
                , Maybe.map editingAudio audioEffect |> Maybe.withDefault Cmd.none
                ]
            )

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
            , editingAudio (Encode.object [ ( "action", Encode.string "stop" ) ])
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

        ChooseFile ->
            ( model, File.Select.file [ "audio/*" ] FilePicked )

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

        ToggleScripture id reference ->
            case findSermon id model.sermons of
                Just sermon ->
                    let
                        options =
                            Api.scriptureOptions sermon

                        current =
                            Dict.get id model.scriptureDrafts
                                |> Maybe.withDefault (Set.fromList sermon.scriptures)

                        updated =
                            if Set.member reference current then
                                Set.remove reference current

                            else
                                Set.insert reference current

                        selected =
                            List.filter (\option -> Set.member option updated) options

                        nextModel =
                            { model
                                | scriptureDrafts = Dict.insert id updated model.scriptureDrafts
                                , scriptureSaveErrors = Set.remove id model.scriptureSaveErrors
                            }
                    in
                    if Set.member id model.scriptureSaving then
                        ( nextModel, Cmd.none )

                    else
                        ( { nextModel | scriptureSaving = Set.insert id model.scriptureSaving }
                        , Api.saveScriptures (ScriptureSaveFinished id selected) id selected
                        )

                Nothing ->
                    ( model, Cmd.none )

        ScriptureSaveFinished id submitted (Ok sermon) ->
            let
                latest =
                    Dict.get id model.scriptureDrafts
                        |> Maybe.map (\selection -> List.filter (\option -> Set.member option selection) (Api.scriptureOptions sermon))
                        |> Maybe.withDefault submitted

                updated =
                    { model
                        | sermons = upsertSermon sermon model.sermons
                        , scriptureSaveErrors = Set.remove id model.scriptureSaveErrors
                    }
            in
            if latest == submitted then
                ( { updated
                    | scriptureDrafts = Dict.remove id updated.scriptureDrafts
                    , scriptureSaving = Set.remove id updated.scriptureSaving
                  }
                , Cmd.none
                )

            else
                ( updated
                , Api.saveScriptures (ScriptureSaveFinished id latest) id latest
                )

        ScriptureSaveFinished id _ (Err _) ->
            ( { model
                | scriptureSaving = Set.remove id model.scriptureSaving
                , scriptureSaveErrors = Set.insert id model.scriptureSaveErrors
              }
            , Cmd.none
            )

        RetryScriptureSave sermon ->
            let
                selected =
                    Dict.get sermon.id model.scriptureDrafts
                        |> Maybe.map (\selection -> List.filter (\option -> Set.member option selection) (Api.scriptureOptions sermon))
                        |> Maybe.withDefault sermon.scriptures
            in
            ( { model
                | scriptureSaving = Set.insert sermon.id model.scriptureSaving
                , scriptureSaveErrors = Set.remove sermon.id model.scriptureSaveErrors
              }
            , Api.saveScriptures (ScriptureSaveFinished sermon.id selected) sermon.id selected
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
        , editingAudioStatus (Editing.AudioStatus >> EditingMsg)
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


findSermon : String -> SermonList -> Maybe Api.Sermon
findSermon id sermonList =
    case sermonList of
        Loaded sermons ->
            List.filter (\sermon -> sermon.id == id) sermons |> List.head

        _ ->
            Nothing


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
