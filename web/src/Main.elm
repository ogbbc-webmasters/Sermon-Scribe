port module Main exposing (main)

import Api
import Browser
import Http
import Json.Decode as Decode
import Set
import Task
import Time
import Types exposing (Model, Msg(..), SermonList(..), UploadState(..))
import View


port pipelineEvents : (Decode.Value -> msg) -> Sub msg


main : Program () Model Msg
main =
    Browser.element
        { init = init
        , update = update
        , view = View.view
        , subscriptions = subscriptions
        }


init : () -> ( Model, Cmd Msg )
init _ =
    ( { sermons = Loading
      , hasPipelineSnapshot = False
      , upload = Idle
      , confirmingDelete = Nothing
      , deleting = Set.empty
      , deletedSermons = Set.empty
      , deleteError = Nothing
      , retrying = Set.empty
      , retryError = Nothing
      , zone = Time.utc
      }
    , Cmd.batch [ Api.fetchSermons GotSermons, Task.perform GotZone Time.here ]
    )



-- UPDATE


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
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
                    ( { model
                        | sermons =
                            Loaded
                                (List.filter
                                    (\sermon -> not (Set.member sermon.id model.deletedSermons))
                                    sermons
                                )
                        , hasPipelineSnapshot = True
                      }
                    , Cmd.none
                    )

                Ok (Api.PipelineUpdate sermon) ->
                    if Set.member sermon.id model.deletedSermons then
                        ( model, Cmd.none )

                    else
                        ( { model | sermons = upsertSermon sermon model.sermons }
                        , Cmd.none
                        )

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

        RetrySermon sermon ->
            ( { model
                | retrying = Set.insert sermon.id model.retrying
                , retryError = Nothing
              }
            , Api.retrySermon (RetryFinished sermon) sermon.id
            )

        RetryFinished original (Ok sermon) ->
            ( { model
                | retrying = Set.remove original.id model.retrying
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
                , retryError = Just "Could not retry. Please try again."
              }
            , Cmd.none
            )

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
