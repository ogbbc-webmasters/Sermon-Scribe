port module Main exposing (main)

import Api
import Browser
import Editor
import Http
import Json.Decode as Decode
import Json.Encode as Encode
import Set
import Task
import Time
import Types exposing (Model, Msg(..), SermonList(..), UploadState(..))
import View


port pipelineEvents : (Decode.Value -> msg) -> Sub msg


port loadTimelineDraft : String -> Cmd msg


port timelineDraftLoaded : (Decode.Value -> msg) -> Sub msg


port saveTimelineDraft : Decode.Value -> Cmd msg


port clearTimelineDraft : String -> Cmd msg


port renderTimeline : Decode.Value -> Cmd msg


port timelineRegionSelected : (Decode.Value -> msg) -> Sub msg


port configurePreview : Decode.Value -> Cmd msg


port previewPlayhead : (Decode.Value -> msg) -> Sub msg


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
      , editing = Nothing
      , hasPipelineSnapshot = False
      , upload = Idle
      , confirmingDelete = Nothing
      , deleting = Set.empty
      , deletedSermons = Set.empty
      , deleteError = Nothing
      , retrying = Set.empty
      , retryError = Nothing
      , rerunning = Set.empty
      , reviewingNormalization = Set.empty
      , normalizationError = Nothing
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
                    let
                        visible =
                            List.filter (\sermon -> not (Set.member sermon.id model.deletedSermons)) sermons

                        ( editing, effect ) =
                            case model.editing of
                                Just editor ->
                                    case List.filter (\sermon -> sermon.id == editor.sermon.id) visible |> List.head of
                                        Just sermon ->
                                            let
                                                ( next, editorEffect ) =
                                                    Editor.pipeline sermon editor
                                            in
                                            ( Just next, editorEffect )

                                        Nothing ->
                                            ( Just editor, Editor.Close )

                                Nothing ->
                                    ( Nothing, Editor.None )

                        updated =
                            { model
                                | sermons =
                                    Loaded visible
                                , hasPipelineSnapshot = True
                                , editing = editing
                            }
                    in
                    performEditor effect updated Cmd.none

                Ok (Api.PipelineUpdate sermon) ->
                    if Set.member sermon.id model.deletedSermons then
                        ( model, Cmd.none )

                    else
                        let
                            ( editing, effect ) =
                                case model.editing of
                                    Just editor ->
                                        let
                                            ( next, editorEffect ) =
                                                Editor.pipeline sermon editor
                                        in
                                        ( Just next, editorEffect )

                                    Nothing ->
                                        ( Nothing, Editor.None )

                            updated =
                                { model | sermons = upsertSermon sermon model.sermons, editing = editing }
                        in
                        performEditor effect updated Cmd.none

                Ok (Api.PipelineDeleted id) ->
                    let
                        closesEditor =
                            model.editing
                                |> Maybe.map (\editor -> editor.sermon.id == id)
                                |> Maybe.withDefault False

                        remainingEditor =
                            case model.editing of
                                Just editor ->
                                    if editor.sermon.id == id then
                                        Nothing

                                    else
                                        model.editing

                                Nothing ->
                                    Nothing
                    in
                    ( { model
                        | deleting = Set.remove id model.deleting
                        , deletedSermons = Set.insert id model.deletedSermons
                        , sermons = removeSermon id model.sermons
                        , editing = remainingEditor
                      }
                    , if closesEditor then
                        Cmd.batch [ configurePreview Encode.null, clearTimelineDraft id ]

                      else
                        Cmd.none
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
            let
                ( editor, command, effect ) =
                    Editor.init sermon

                updated =
                    { model
                        | sermons = upsertSermon sermon model.sermons
                        , editing = Just editor
                        , reviewingNormalization = Set.remove sermon.id model.reviewingNormalization
                    }
            in
            performEditor effect updated (Cmd.map EditorMsg command)

        NormalizationReviewed original (Err _) ->
            ( { model
                | reviewingNormalization = Set.remove original.id model.reviewingNormalization
                , normalizationError = Just "Could not save the normalization review. Please try again."
              }
            , Cmd.none
            )

        OpenEditor sermon ->
            let
                ( editor, command, effect ) =
                    Editor.init sermon
            in
            performEditor effect { model | editing = Just editor } (Cmd.map EditorMsg command)

        EditorMsg editorMsg ->
            case model.editing of
                Just editor ->
                    let
                        ( next, command, effect ) =
                            Editor.update editorMsg editor
                    in
                    performEditor effect { model | editing = Just next } (Cmd.map EditorMsg command)

                Nothing ->
                    ( model, Cmd.none )

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
        , timelineDraftLoaded (EditorMsg << Editor.GotDraft)
        , timelineRegionSelected (EditorMsg << Editor.CanvasSelect)
        , previewPlayhead (EditorMsg << Editor.Playhead)
        , case model.upload of
            Uploading _ ->
                Http.track Api.uploadTracker UploadProgress

            _ ->
                Sub.none
        ]


performEditor : Editor.Effect -> Model -> Cmd Msg -> ( Model, Cmd Msg )
performEditor effect model command =
    case effect of
        Editor.None ->
            ( model, command )

        Editor.LoadDraft id ->
            ( model, Cmd.batch [ command, loadTimelineDraft id ] )

        Editor.SaveDraft id value render preview ->
            ( model, Cmd.batch [ command, saveTimelineDraft (Encode.object [ ( "id", Encode.string id ), ( "draft", value ) ]), renderTimeline render, configurePreview preview ] )

        Editor.ClearDraft id ->
            ( model, Cmd.batch [ command, clearTimelineDraft id ] )

        Editor.Render value preview ->
            ( model, Cmd.batch [ command, renderTimeline value, configurePreview preview ] )

        Editor.Complete id value preview ->
            ( model, Cmd.batch [ command, renderTimeline value, configurePreview preview, clearTimelineDraft id ] )

        Editor.Preview value ->
            ( model, Cmd.batch [ command, configurePreview value, clearTimelineDraft (model.editing |> Maybe.map (.sermon >> .id) |> Maybe.withDefault "") ] )

        Editor.Close ->
            ( { model | editing = Nothing }, Cmd.batch [ command, configurePreview Encode.null ] )

        Editor.ApprovedSermon sermon ->
            ( { model | editing = Nothing, sermons = upsertSermon sermon model.sermons }, Cmd.batch [ command, clearTimelineDraft sermon.id, configurePreview Encode.null ] )


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
